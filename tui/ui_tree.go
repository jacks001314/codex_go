package tui

// Rust parity: codex-rs/tui/src/ui_tree.rs and ui_tree/render.rs (#57970).
//
// A bounded, display-only subset of the `codex-tui/1` tree contract. Producers
// normalize text into leaf nodes and arrays into composite nodes; unsupported
// primitives and properties fail deserialization. This slice renders one
// horizontal line of plain text, links, rows, spacers, dividers, and numeric
// gaps. Links underline their text and carry an explicit hyperlink range so the
// footer can truncate without moving the destination onto other cells.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

const (
	// UITreeMaxDepth bounds nested composite nodes.
	UITreeMaxDepth = 16
	// UITreeMaxNodes bounds the total nodes visited in one render.
	UITreeMaxNodes = 256
	// UITreeMaxTextBytes bounds the text bytes visited in one render.
	UITreeMaxTextBytes = 16 * 1024
	// UITreeMaxGap clamps a row gap in points before conversion to cells.
	UITreeMaxGap = 32.0
)

// UINodeKind identifies the serialized node type.
type UINodeKind string

const (
	UINodeText    UINodeKind = "text"
	UINodeLink    UINodeKind = "link"
	UINodeRow     UINodeKind = "row"
	UINodeSpacer  UINodeKind = "spacer"
	UINodeDivider UINodeKind = "divider"
)

// LinkHref is an absolute HTTP(S) destination safe to embed in a terminal
// hyperlink.
type LinkHref struct {
	value string
}

// NewLinkHref validates an absolute HTTP(S) URL without control characters.
func NewLinkHref(raw string) (LinkHref, error) {
	destination, ok := WebDestination(raw)
	if !ok || destination != raw {
		return LinkHref{}, errors.New("link href must be an absolute HTTP(S) URL without control characters")
	}
	return LinkHref{value: raw}, nil
}

func (l LinkHref) String() string { return l.value }

func (l LinkHref) MarshalJSON() ([]byte, error) { return json.Marshal(l.value) }

func (l *LinkHref) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	href, err := NewLinkHref(raw)
	if err != nil {
		return err
	}
	*l = href
	return nil
}

// UINode is one display node. Children entries may be nil (serialized null).
type UINode struct {
	Kind     UINodeKind
	Key      json.RawMessage
	Text     string
	Href     LinkHref
	Title    *string
	Gap      *float64
	Children []*UINode
}

// ParseUITree decodes a JSON tree. A JSON null decodes to a nil tree.
func ParseUITree(data []byte) (*UINode, error) {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	return decodeUINode(trimmed)
}

// MustUITree is ParseUITree for fixtures that are expected to decode.
func MustUITree(data []byte) *UINode {
	node, err := ParseUITree(data)
	if err != nil {
		panic(err)
	}
	return node
}

func decodeUINode(data []byte) (*UINode, error) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	var kind string
	if raw, ok := fields["type"]; !ok {
		return nil, errors.New("ui node missing type")
	} else if err := json.Unmarshal(raw, &kind); err != nil {
		return nil, err
	}
	node := &UINode{Kind: UINodeKind(kind)}
	if raw, ok := fields["key"]; ok {
		node.Key = append(json.RawMessage(nil), raw...)
	}
	var props map[string]json.RawMessage
	if raw, ok := fields["props"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		props = map[string]json.RawMessage{}
		if err := json.Unmarshal(raw, &props); err != nil {
			return nil, err
		}
	}
	var childrenRaw json.RawMessage
	if raw, ok := fields["children"]; ok {
		childrenRaw = raw
	}
	allowed := map[string]bool{"type": true, "key": true, "props": true, "children": true}
	for name := range fields {
		if !allowed[name] {
			return nil, fmt.Errorf("ui node field %q is not supported", name)
		}
	}
	switch node.Kind {
	case UINodeText:
		if childrenRaw != nil {
			return nil, errors.New("ui text node must not carry children")
		}
		raw, ok := props["text"]
		if !ok {
			return nil, errors.New("ui text node missing props.text")
		}
		if err := rejectUnknownProps(props, "text"); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &node.Text); err != nil {
			return nil, err
		}
	case UINodeLink:
		raw, ok := props["href"]
		if !ok {
			return nil, errors.New("ui link node missing props.href")
		}
		if err := json.Unmarshal(raw, &node.Href); err != nil {
			return nil, err
		}
		if rawTitle, ok := props["title"]; ok && !bytes.Equal(bytes.TrimSpace(rawTitle), []byte("null")) {
			var title string
			if err := json.Unmarshal(rawTitle, &title); err != nil {
				return nil, err
			}
			node.Title = &title
		}
		if err := rejectUnknownProps(props, "href", "title"); err != nil {
			return nil, err
		}
		children, err := decodeChildren(childrenRaw)
		if err != nil {
			return nil, err
		}
		node.Children = children
	case UINodeRow:
		if rawGap, ok := props["gap"]; ok && !bytes.Equal(bytes.TrimSpace(rawGap), []byte("null")) {
			var gap float64
			if err := json.Unmarshal(rawGap, &gap); err != nil {
				return nil, err
			}
			node.Gap = &gap
		}
		if err := rejectUnknownProps(props, "gap"); err != nil {
			return nil, err
		}
		children, err := decodeChildren(childrenRaw)
		if err != nil {
			return nil, err
		}
		node.Children = children
	case UINodeSpacer, UINodeDivider:
		if childrenRaw != nil {
			return nil, errors.New("ui leaf node must not carry children")
		}
		if len(props) > 0 {
			return nil, errors.New("ui leaf node must not carry props")
		}
	default:
		return nil, fmt.Errorf("unsupported ui node type %q", kind)
	}
	return node, nil
}

func rejectUnknownProps(props map[string]json.RawMessage, allowed ...string) error {
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}
	for name := range props {
		if !allowedSet[name] {
			return fmt.Errorf("ui node prop %q is not supported", name)
		}
	}
	return nil
}

func decodeChildren(raw json.RawMessage) ([]*UINode, error) {
	if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	children := make([]*UINode, 0, len(entries))
	for _, entry := range entries {
		child, err := decodeUINode(entry)
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	return children, nil
}

// MarshalJSON serializes the node in the `codex-tui/1` shape.
func (n *UINode) MarshalJSON() ([]byte, error) {
	if n == nil {
		return []byte("null"), nil
	}
	out := map[string]json.RawMessage{}
	out["type"], _ = json.Marshal(string(n.Kind))
	if len(n.Key) > 0 {
		out["key"] = n.Key
	}
	switch n.Kind {
	case UINodeText:
		props, _ := json.Marshal(map[string]any{"text": n.Text})
		out["props"] = props
	case UINodeLink:
		props := map[string]any{"href": n.Href.String()}
		if n.Title != nil {
			props["title"] = *n.Title
		}
		encodedProps, _ := json.Marshal(props)
		out["props"] = encodedProps
		children, err := marshalChildren(n.Children)
		if err != nil {
			return nil, err
		}
		out["children"] = children
	case UINodeRow:
		props := map[string]any{}
		if n.Gap != nil {
			props["gap"] = *n.Gap
		}
		encodedProps, _ := json.Marshal(props)
		out["props"] = encodedProps
		children, err := marshalChildren(n.Children)
		if err != nil {
			return nil, err
		}
		out["children"] = children
	}
	return json.Marshal(out)
}

func marshalChildren(children []*UINode) (json.RawMessage, error) {
	encoded := make([]json.RawMessage, 0, len(children))
	for _, child := range children {
		raw, err := child.MarshalJSON()
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, raw)
	}
	return json.Marshal(encoded)
}

// UILine is one rendered display row: plain text plus explicit hyperlink
// column ranges.
type UILine struct {
	Text       string
	Hyperlinks []TerminalHyperlink
}

// Width returns the display width of the rendered text.
func (l UILine) Width() int { return DisplayWidth(l.Text) }

// Render emits the row with OSC-8 hyperlinks applied to their exact columns.
func (l UILine) Render() string {
	if len(l.Hyperlinks) == 0 || l.Text == "" {
		return l.Text
	}
	ranges := append([]TerminalHyperlink(nil), l.Hyperlinks...)
	sort.SliceStable(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	var out strings.Builder
	column := 0
	active := ""
	graphemes := uniseg.NewGraphemes(l.Text)
	for graphemes.Next() {
		grapheme := graphemes.Str()
		start, _ := graphemes.Positions()
		destination := ""
		for _, link := range ranges {
			if column >= link.Start && column < link.End {
				destination = link.Destination
				break
			}
		}
		if destination != active {
			if active != "" {
				out.WriteString(osc8CloseSequence)
			}
			if destination != "" {
				out.WriteString(osc8OpenSequence(destination))
			}
			active = destination
		}
		out.WriteString(l.Text[start : start+len(grapheme)])
		column += DisplayWidth(grapheme)
	}
	if active != "" {
		out.WriteString(osc8CloseSequence)
	}
	return out.String()
}

const osc8CloseSequence = "\x1b]8;;\x07"

func osc8OpenSequence(destination string) string {
	safe, ok := WebDestination(destination)
	if !ok {
		return ""
	}
	return "\x1b]8;;" + safe + "\x07"
}

// RenderLine renders the visible prefix of the tree into one display row. On
// overflow it fits an unlinked ellipsis inside the supplied width.
func (n *UINode) RenderLine(width int) UILine {
	if n == nil || width <= 0 {
		return UILine{}
	}
	budget := &uiRenderBudget{nodes: UITreeMaxNodes, textBytes: UITreeMaxTextBytes}
	line := uiNodeLine(n, 0, budget)
	if line == nil {
		return UILine{}
	}
	if DisplayWidth(line.Text) > width {
		keep := width - 1
		line.Text = TruncateToWidth(line.Text, keep) + "…"
		line.Hyperlinks = ClipHyperlinksToWidth(line.Hyperlinks, keep)
	}
	return *line
}

type uiRenderBudget struct {
	nodes     int
	textBytes int
}

func uiNodeLine(node *UINode, depth int, budget *uiRenderBudget) *UILine {
	if node == nil || budget.nodes == 0 {
		return nil
	}
	budget.nodes--
	if depth > UITreeMaxDepth {
		return nil
	}
	switch node.Kind {
	case UINodeText:
		raw := node.Text
		if budget.textBytes == 0 && raw != "" {
			return nil
		}
		end := len(raw)
		if end > budget.textBytes {
			end = budget.textBytes
		}
		for end > 0 && end < len(raw) && !utf8.RuneStart(raw[end]) {
			end--
		}
		first := strings.SplitN(raw[:end], "\n", 2)[0]
		budget.textBytes -= len(first)
		visible := strings.ReplaceAll(sanitizeUITreeText(first), "\t", "    ")
		return &UILine{Text: visible}
	case UINodeLink:
		line := uiRowLine(node.Children, 0, depth, budget)
		if line == nil {
			line = &UILine{}
		}
		if width := DisplayWidth(line.Text); width > 0 {
			line.Hyperlinks = []TerminalHyperlink{{Start: 0, End: width, Destination: node.Href.String()}}
		}
		return line
	case UINodeRow:
		gap := 0
		if node.Gap != nil {
			points := math.Max(0, math.Min(UITreeMaxGap, *node.Gap))
			gap = int(math.Ceil(points / 2))
		}
		return uiRowLine(node.Children, gap, depth, budget)
	case UINodeSpacer:
		return &UILine{Text: " "}
	case UINodeDivider:
		return &UILine{Text: "│"}
	}
	return nil
}

func uiRowLine(children []*UINode, gap int, depth int, budget *uiRenderBudget) *UILine {
	var result *UILine
	used := 0
	for _, child := range children {
		if budget.nodes == 0 {
			break
		}
		childLine := uiNodeLine(child, depth+1, budget)
		if childLine == nil {
			continue
		}
		spacing := 0
		if result != nil {
			spacing = gap
		} else {
			result = &UILine{}
		}
		if spacing > 0 {
			result.Text += strings.Repeat(" ", spacing)
		}
		offset := used + spacing
		used = offset + DisplayWidth(childLine.Text)
		result.Text += childLine.Text
		for _, link := range childLine.Hyperlinks {
			result.Hyperlinks = append(result.Hyperlinks, TerminalHyperlink{
				Start:       link.Start + offset,
				End:         link.End + offset,
				Destination: link.Destination,
			})
		}
	}
	return result
}

// ClipHyperlinksToWidth clamps ranges to width and drops empty ones.
func ClipHyperlinksToWidth(links []TerminalHyperlink, width int) []TerminalHyperlink {
	if len(links) == 0 {
		return nil
	}
	clipped := make([]TerminalHyperlink, 0, len(links))
	for _, link := range links {
		start := link.Start
		end := link.End
		if start > width {
			start = width
		}
		if end > width {
			end = width
		}
		if end <= start {
			continue
		}
		clipped = append(clipped, TerminalHyperlink{Start: start, End: end, Destination: link.Destination})
	}
	if len(clipped) == 0 {
		return nil
	}
	return clipped
}

// sanitizeUITreeText mirrors history_cell.SanitizeUserText without importing
// that package (which itself imports tui). It removes CSI escapes and control
// characters while preserving tabs and newlines.
func sanitizeUITreeText(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	for index := 0; index < len(text); {
		r, size := utf8.DecodeRuneInString(text[index:])
		if r == 0x1b {
			rest := text[index+size:]
			if strings.HasPrefix(rest, "[") {
				if end := csiUITreeEnd(rest); end >= 0 {
					index += size + end
					continue
				}
				return out.String()
			}
			index += size
			continue
		}
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			index += size
			continue
		}
		out.WriteString(text[index : index+size])
		index += size
	}
	return out.String()
}

func csiUITreeEnd(rest string) int {
	for offset := 1; offset < len(rest); offset++ {
		if final := rest[offset]; final >= '@' && final <= '~' {
			return offset + 1
		}
	}
	return -1
}
