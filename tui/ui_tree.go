package tui

// Rust parity: codex-rs/tui/src/ui_tree.rs, ui_tree/decode.rs and
// ui_tree/render.rs (#57970, #59518).
//
// The wire tree is a display-only subset of the `codex-tui/1` contract. A
// whole-tree decode validates JSON size, depth, node count and text bytes so
// rendering never silently drops content outside the viewport. Text is a string
// leaf or a `text` element with children; rows carry an explicit gap in terminal
// columns. Links underline their text and carry an explicit hyperlink range.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

const (
	// UITreeMaxJSONBytes bounds the complete wire JSON before parsing.
	UITreeMaxJSONBytes = 64 * 1024
	// UITreeMaxDepth bounds nested composite nodes.
	UITreeMaxDepth = 16
	// UITreeMaxNodes bounds non-null nodes in a decoded tree.
	UITreeMaxNodes = 256
	// UITreeMaxTextBytes bounds the total UTF-8 content of string leaves.
	UITreeMaxTextBytes = 16 * 1024
	// UITreeMaxGap is the largest JavaScript-safe non-negative row gap.
	UITreeMaxGap uint64 = (1 << 53) - 1
	// UITreeMaxLinkBytes bounds a terminal hyperlink destination.
	UITreeMaxLinkBytes = 8 * 1024
)

// UINodeKind identifies the serialized node type.
type UINodeKind string

const (
	// UINodeText is a text element containing child nodes.
	UINodeText UINodeKind = "text"
	// UINodeLink is a hyperlink containing child nodes.
	UINodeLink UINodeKind = "link"
	// UINodeRow lays its children out horizontally.
	UINodeRow UINodeKind = "row"
	// UINodeString is a bare JSON string leaf.
	UINodeString UINodeKind = "string"
)

// LinkHref is an absolute HTTP(S) destination safe to embed in a terminal
// hyperlink.
type LinkHref struct {
	value string
}

// NewLinkHref validates an absolute HTTP(S) URL without control characters and
// at most 8 KiB in UTF-8.
func NewLinkHref(raw string) (LinkHref, error) {
	destination, ok := WebDestination(raw)
	if !ok || destination != raw || len(raw) > UITreeMaxLinkBytes {
		return LinkHref{}, errors.New("link href must be an absolute HTTP(S) URL of at most 8 KiB without control characters")
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
// For UINodeString, Text carries the JSON string leaf. For UINodeText, Children
// carry all content. Gap is the explicit row spacing in terminal columns.
type UINode struct {
	Kind     UINodeKind
	Text     string
	Href     LinkHref
	Title    *string
	Gap      uint64
	Children []*UINode
}

// ParseUITree decodes one whole JSON tree and validates every node, including
// content outside the eventual viewport. A JSON null decodes to a nil tree.
func ParseUITree(data []byte) (*UINode, error) {
	if len(data) > UITreeMaxJSONBytes {
		return nil, errors.New("UI tree JSON exceeds 64 KiB")
	}
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	node, err := decodeUINode(trimmed)
	if err != nil {
		return nil, err
	}
	if err := validateUITree(node); err != nil {
		return nil, err
	}
	return node, nil
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
	trimmed := bytes.TrimSpace(data)
	switch {
	case len(trimmed) == 0:
		return nil, errors.New("empty ui node")
	case bytes.Equal(trimmed, []byte("null")):
		return nil, nil
	case trimmed[0] == '"':
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, err
		}
		return &UINode{Kind: UINodeString, Text: text}, nil
	case trimmed[0] != '{':
		return nil, errors.New("ui node must be an object, string, or null")
	}
	fields, err := decodeJSONObject(trimmed)
	if err != nil {
		return nil, err
	}
	for name := range fields {
		switch name {
		case "type", "props", "children":
		default:
			return nil, fmt.Errorf("ui node field %q is not supported", name)
		}
	}
	var kind string
	if raw, ok := fields["type"]; !ok {
		return nil, errors.New("ui node missing type")
	} else if err := json.Unmarshal(raw, &kind); err != nil {
		return nil, err
	}
	node := &UINode{Kind: UINodeKind(kind)}
	props, err := decodeUIProps(fields["props"])
	if err != nil {
		return nil, err
	}
	childrenRaw, hasChildren := fields["children"]
	switch node.Kind {
	case UINodeText:
		if len(props) != 0 {
			return nil, errors.New("ui text node props must be empty")
		}
		children, err := decodeRequiredUIChildren(childrenRaw, hasChildren)
		if err != nil {
			return nil, err
		}
		node.Children = children
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
		children, err := decodeRequiredUIChildren(childrenRaw, hasChildren)
		if err != nil {
			return nil, err
		}
		node.Children = children
	case UINodeRow:
		raw, ok := props["gap"]
		if !ok {
			return nil, errors.New("ui row node missing props.gap")
		}
		gap, err := decodeUIRowGap(raw)
		if err != nil {
			return nil, err
		}
		node.Gap = gap
		if err := rejectUnknownProps(props, "gap"); err != nil {
			return nil, err
		}
		children, err := decodeRequiredUIChildren(childrenRaw, hasChildren)
		if err != nil {
			return nil, err
		}
		node.Children = children
	default:
		return nil, fmt.Errorf("unsupported ui node type %q", kind)
	}
	return node, nil
}

func decodeUIProps(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if raw == nil {
		return map[string]json.RawMessage{}, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errors.New("ui node props must be an object")
	}
	return decodeJSONObject(raw)
}

// decodeJSONObject rejects duplicate keys, which encoding/json's map decoder
// would otherwise collapse before the tree contract can inspect them.
func decodeJSONObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, errors.New("ui node must be an object")
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, errors.New("ui object key must be a string")
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate ui object field %q", name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = append(json.RawMessage(nil), value...)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("unexpected trailing ui JSON")
		}
		return nil, err
	}
	return fields, nil
}

func decodeRequiredUIChildren(raw json.RawMessage, present bool) ([]*UINode, error) {
	if !present {
		return nil, errors.New("ui composite node missing children")
	}
	return decodeUIChildren(raw)
}

func decodeUIRowGap(raw json.RawMessage) (uint64, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, err
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, errors.New("ui row gap must be a non-negative integer")
	}
	text := number.String()
	if gap, err := strconv.ParseUint(text, 10, 64); err == nil {
		if gap > UITreeMaxGap {
			return 0, errors.New("ui row gap exceeds the maximum safe integer")
		}
		return gap, nil
	}
	gap, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(gap) || math.IsInf(gap, 0) || gap < 0 || math.Trunc(gap) != gap || gap > float64(UITreeMaxGap) {
		return 0, errors.New("ui row gap must be a non-negative JavaScript-safe integer")
	}
	return uint64(gap), nil
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

func decodeUIChildren(raw json.RawMessage) ([]*UINode, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errors.New("ui node children must be an array")
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

func validateUITree(root *UINode) error {
	if root == nil {
		return nil
	}
	type pendingNode struct {
		node  *UINode
		depth int
	}
	pending := []pendingNode{{node: root}}
	nodes := 0
	textBytes := 0
	for len(pending) > 0 {
		entry := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if entry.depth > UITreeMaxDepth {
			return errors.New("UI tree exceeds maximum depth 16")
		}
		nodes++
		if nodes > UITreeMaxNodes {
			return errors.New("UI tree exceeds 256 non-null nodes")
		}
		if entry.node.Kind == UINodeString {
			textBytes += len(entry.node.Text)
			if textBytes > UITreeMaxTextBytes {
				return errors.New("UI tree exceeds 16 KiB of text")
			}
			continue
		}
		for index := len(entry.node.Children) - 1; index >= 0; index-- {
			if child := entry.node.Children[index]; child != nil {
				pending = append(pending, pendingNode{node: child, depth: entry.depth + 1})
			}
		}
	}
	return nil
}

// MarshalJSON serializes the node in the `codex-tui/1` shape.
func (n *UINode) MarshalJSON() ([]byte, error) {
	if n == nil {
		return []byte("null"), nil
	}
	if n.Kind == UINodeString {
		return json.Marshal(n.Text)
	}
	out := map[string]json.RawMessage{}
	out["type"], _ = json.Marshal(string(n.Kind))
	switch n.Kind {
	case UINodeText:
		children, err := marshalUIChildren(n.Children)
		if err != nil {
			return nil, err
		}
		out["children"] = children
	case UINodeLink:
		props := map[string]any{"href": n.Href.String()}
		if n.Title != nil {
			props["title"] = *n.Title
		}
		encodedProps, _ := json.Marshal(props)
		out["props"] = encodedProps
		children, err := marshalUIChildren(n.Children)
		if err != nil {
			return nil, err
		}
		out["children"] = children
	case UINodeRow:
		encodedProps, _ := json.Marshal(map[string]any{"gap": n.Gap})
		out["props"] = encodedProps
		children, err := marshalUIChildren(n.Children)
		if err != nil {
			return nil, err
		}
		out["children"] = children
	default:
		return nil, fmt.Errorf("unsupported ui node type %q", n.Kind)
	}
	return json.Marshal(out)
}

func marshalUIChildren(children []*UINode) (json.RawMessage, error) {
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
	line := uiNodeLine(n, width)
	if line.Width() > width {
		keep := width - 1
		line.Text = TruncateToWidth(line.Text, keep) + "…"
		line.Hyperlinks = ClipHyperlinksToWidth(line.Hyperlinks, keep)
	}
	return line
}

func uiNodeLine(node *UINode, width int) UILine {
	if node == nil {
		return UILine{}
	}
	switch node.Kind {
	case UINodeString:
		raw := boundedUITreeText(node.Text)
		first := strings.SplitN(raw, "\n", 2)[0]
		return UILine{Text: strings.ReplaceAll(sanitizeUITreeText(first), "\t", "    ")}
	case UINodeText:
		return uiRowLine(node.Children, 0, width)
	case UINodeLink:
		line := uiRowLine(node.Children, 0, width)
		if lineWidth := line.Width(); lineWidth > 0 {
			line.Hyperlinks = []TerminalHyperlink{{Start: 0, End: lineWidth, Destination: node.Href.String()}}
		}
		return line
	case UINodeRow:
		return uiRowLine(node.Children, node.Gap, width)
	default:
		return UILine{}
	}
}

func boundedUITreeText(raw string) string {
	if len(raw) <= UITreeMaxTextBytes {
		return raw
	}
	end := UITreeMaxTextBytes
	for end > 0 && end < len(raw) && !utf8.RuneStart(raw[end]) {
		end--
	}
	return raw[:end]
}

func uiRowLine(children []*UINode, gap uint64, width int) UILine {
	var result UILine
	used := 0
	for _, child := range children {
		childLine := uiNodeLine(child, width)
		childWidth := childLine.Width()
		if childWidth == 0 {
			continue
		}
		if used > 0 && gap > 0 {
			// One column past the viewport is sufficient to detect overflow.
			available := width + 1 - used
			if available < 0 {
				available = 0
			}
			spacing := gap
			if spacing > uint64(available) {
				spacing = uint64(available)
			}
			if spacing > 0 {
				result.Text += strings.Repeat(" ", int(spacing))
				used += int(spacing)
			}
		}
		offset := used
		result.Text += childLine.Text
		for _, link := range childLine.Hyperlinks {
			result.Hyperlinks = append(result.Hyperlinks, TerminalHyperlink{
				Start:       link.Start + offset,
				End:         link.End + offset,
				Destination: link.Destination,
			})
		}
		used += childWidth
		if used > width {
			break
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
