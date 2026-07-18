package status

import (
	"strings"

	codextui "codex_go/tui"
)

func Join(left string, right string) string {
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	return left + " | " + right
}

type FieldFormatter struct {
	Indent      string
	LabelWidth  int
	ValueOffset int
	ValueIndent string
}

const FieldFormatterIndent = " "

func NewFieldFormatter(labels []string) FieldFormatter {
	labelWidth := 0
	for _, label := range labels {
		if width := codextui.DisplayWidth(label); width > labelWidth {
			labelWidth = width
		}
	}
	valueOffset := codextui.DisplayWidth(FieldFormatterIndent) + labelWidth + 1 + 3
	return FieldFormatter{
		Indent:      FieldFormatterIndent,
		LabelWidth:  labelWidth,
		ValueOffset: valueOffset,
		ValueIndent: strings.Repeat(" ", valueOffset),
	}
}

func (f FieldFormatter) Line(label string, value string) string {
	return f.LabelPrefix(label) + value
}

func (f FieldFormatter) Continuation(value string) string {
	return f.ValueIndent + value
}

func (f FieldFormatter) ValueWidth(availableInnerWidth int) int {
	if availableInnerWidth <= f.ValueOffset {
		return 0
	}
	return availableInnerWidth - f.ValueOffset
}

func (f FieldFormatter) LabelPrefix(label string) string {
	labelWidth := codextui.DisplayWidth(label)
	padding := 3
	if f.LabelWidth > labelWidth {
		padding += f.LabelWidth - labelWidth
	}
	return f.Indent + label + ":" + strings.Repeat(" ", padding)
}

func PushLabel(labels *[]string, seen map[string]struct{}, label string) {
	if labels == nil || seen == nil {
		return
	}
	if _, ok := seen[label]; ok {
		return
	}
	seen[label] = struct{}{}
	*labels = append(*labels, label)
}

func LineDisplayWidth(line string) int {
	return codextui.DisplayWidth(line)
}

func TruncateLineToWidth(line string, maxWidth int) string {
	return codextui.TruncateToWidth(line, maxWidth)
}
