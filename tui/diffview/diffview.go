// Package diffview provides a reusable terminal diff viewer component.
// It supports unified and split (side-by-side) layouts with optional
// syntax highlighting and line numbering.
package diffview

import (
	"strings"

	codextui "codex_go/tui"
	"github.com/charmbracelet/lipgloss"
)

// Layout controls how the diff is displayed.
type Layout int

const (
	// LayoutUnified shows a single column with + and - prefixed lines.
	LayoutUnified Layout = iota

	// LayoutSplit shows two side-by-side columns: old on the left, new on the right.
	LayoutSplit
)

// LineKind classifies a single line in a diff hunk.
type LineKind int

const (
	LineContext LineKind = iota
	LineInsert
	LineDelete
)

// DiffLine represents a single line in the diff output.
type DiffLine struct {
	Kind    LineKind
	Content string
	OldNum  int // line number in old file, 0 if not applicable
	NewNum  int // line number in new file, 0 if not applicable
}

// Hunk represents one contiguous change block.
type Hunk struct {
	OldStart int
	NewStart int
	Lines    []DiffLine
}

// FileDiff represents the diff for a single file.
type FileDiff struct {
	Path    string
	OldPath string // set if file was renamed
	Hunks   []Hunk
}

// View renders a structured diff into terminal output.
// It wraps a set of FileDiffs and renders them with the given configuration.
type View struct {
	Files        []FileDiff
	Layout       Layout
	ShowLineNum  bool
	ContextLines int
	Width        int

	// Styles
	AddFg     lipgloss.Color
	AddBg     lipgloss.Color
	DelFg     lipgloss.Color
	DelBg     lipgloss.Color
	ContextFg lipgloss.Color
	HeaderFg  lipgloss.Color

	maxLineNumWidth int
}

// NewView creates a diff view with sensible default styles.
func NewView(width int) *View {
	return &View{
		Layout:       LayoutUnified,
		ShowLineNum:  true,
		ContextLines: 3,
		Width:        width,
		AddFg:        lipgloss.Color("2"),  // green
		AddBg:        lipgloss.Color("22"), // dark green
		DelFg:        lipgloss.Color("1"),  // red
		DelBg:        lipgloss.Color("52"), // dark red
		ContextFg:    lipgloss.Color("8"),  // dim
		HeaderFg:     lipgloss.Color("12"), // blue
	}
}

// AddFile appends a file diff to the view.
func (v *View) AddFile(fd FileDiff) {
	v.Files = append(v.Files, fd)
}

// Render produces the full terminal output string.
func (v *View) Render() string {
	if v.Width <= 0 {
		v.Width = 80
	}
	v.computeMaxLineNumWidth()

	var sections []string
	for i, fd := range v.Files {
		if i > 0 {
			sections = append(sections, "")
		}
		sections = append(sections, v.renderFileHeader(fd))
		for _, h := range fd.Hunks {
			sections = append(sections, v.renderHunk(h))
		}
	}
	return strings.Join(sections, "\n")
}

func (v *View) renderFileHeader(fd FileDiff) string {
	header := "diff"
	if fd.OldPath != "" {
		header += " --git a/" + fd.OldPath + " b/" + fd.Path
	} else {
		header += " " + fd.Path
	}
	return lipgloss.NewStyle().Foreground(v.HeaderFg).Bold(true).Render(header)
}

func (v *View) renderHunk(h Hunk) string {
	switch v.Layout {
	case LayoutSplit:
		return v.renderHunkSplit(h)
	default:
		return v.renderHunkUnified(h)
	}
}

func (v *View) renderHunkUnified(h Hunk) string {
	var lines []string
	for _, dl := range h.Lines {
		var prefix string
		var fg lipgloss.Color
		var bg lipgloss.Color

		switch dl.Kind {
		case LineInsert:
			prefix = "+"
			fg = v.AddFg
			bg = v.AddBg
		case LineDelete:
			prefix = "-"
			fg = v.DelFg
			bg = v.DelBg
		default:
			prefix = " "
			fg = v.ContextFg
		}

		lineNum := ""
		if v.ShowLineNum {
			lineNum = v.formatUnifiedLineNum(dl)
		}
		content := v.truncateLine(lineNum + prefix + dl.Content)
		styled := lipgloss.NewStyle().Foreground(fg).Background(bg).Render(content)
		lines = append(lines, styled)
	}
	return strings.Join(lines, "\n")
}

func (v *View) renderHunkSplit(h Hunk) string {
	// For split view, render old (left) and new (right) side-by-side
	halfWidth := (v.Width - 1) / 2 // -1 for separator

	var lines []string
	for _, dl := range h.Lines {
		oldLine := ""
		newLine := ""

		switch dl.Kind {
		case LineInsert:
			newLine = "+" + dl.Content
		case LineDelete:
			oldLine = "-" + dl.Content
		default:
			oldLine = " " + dl.Content
			newLine = " " + dl.Content
		}

		oldStyled := v.padOrTruncate(oldLine, halfWidth, v.DelFg, v.DelBg)
		newStyled := v.padOrTruncate(newLine, halfWidth, v.AddFg, v.AddBg)
		separator := lipgloss.NewStyle().Foreground(v.ContextFg).Render("│")
		lines = append(lines, oldStyled+separator+newStyled)
	}
	return strings.Join(lines, "\n")
}

func (v *View) formatUnifiedLineNum(dl DiffLine) string {
	if dl.OldNum > 0 && dl.NewNum > 0 {
		// Context line: show first number
		return v.padLineNum(dl.OldNum) + " "
	}
	if dl.OldNum > 0 {
		return v.padLineNum(dl.OldNum) + " "
	}
	if dl.NewNum > 0 {
		return strings.Repeat(" ", v.maxLineNumWidth) + " "
	}
	return strings.Repeat(" ", v.maxLineNumWidth) + " "
}

func (v *View) padLineNum(n int) string {
	s := itoa(n)
	pad := v.maxLineNumWidth - len(s)
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + s
}

func (v *View) truncateLine(line string) string {
	if v.Width <= 0 || codextui.DisplayWidth(line) <= v.Width {
		return line
	}
	if v.Width <= 3 {
		return codextui.TruncateToWidth(line, v.Width)
	}
	return codextui.TruncateWithEllipsis(line, v.Width)
}

func (v *View) padOrTruncate(line string, width int, fg, bg lipgloss.Color) string {
	lineWidth := codextui.DisplayWidth(line)
	if lineWidth > width {
		line = codextui.TruncateWithEllipsis(line, width)
	} else {
		line = line + strings.Repeat(" ", width-lineWidth)
	}
	return lipgloss.NewStyle().Foreground(fg).Background(bg).Render(line)
}

func (v *View) computeMaxLineNumWidth() {
	maxNum := 0
	for _, fd := range v.Files {
		for _, h := range fd.Hunks {
			for _, dl := range h.Lines {
				if dl.OldNum > maxNum {
					maxNum = dl.OldNum
				}
				if dl.NewNum > maxNum {
					maxNum = dl.NewNum
				}
			}
		}
	}
	v.maxLineNumWidth = len(itoa(maxNum))
	if v.maxLineNumWidth < 4 {
		v.maxLineNumWidth = 4
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// ParseUnifiedDiff parses a standard unified diff string into FileDiffs.
// This is a minimal parser that handles common cases.
func ParseUnifiedDiff(diffText string) []FileDiff {
	if strings.TrimSpace(diffText) == "" {
		return nil
	}

	var files []FileDiff
	var current *FileDiff
	var currentHunk *Hunk

	lines := strings.Split(diffText, "\n")
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")

		switch {
		case strings.HasPrefix(line, "diff --git "):
			if current != nil {
				files = append(files, *current)
			}
			current = &FileDiff{}
			currentHunk = nil
			parts := strings.Fields(line)
			for _, p := range parts {
				if strings.HasPrefix(p, "a/") && strings.HasPrefix(parts[len(parts)-1], "b/") {
					current.OldPath = strings.TrimPrefix(p, "a/")
				}
				if strings.HasPrefix(p, "b/") {
					current.Path = strings.TrimPrefix(p, "b/")
				}
			}
			if current.Path == "" && current.OldPath != "" {
				current.Path = current.OldPath
			}

		case strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- "):
			if current != nil && current.Path == "" {
				path := strings.TrimPrefix(line, "+++ ")
				path = strings.TrimPrefix(path, "b/")
				current.Path = path
			}

		case strings.HasPrefix(line, "@@ "):
			if current == nil {
				current = &FileDiff{Path: "unknown"}
			}
			oldStart, newStart := parseHunkHeader(line)
			currentHunk = &Hunk{OldStart: oldStart, NewStart: newStart}
			current.Hunks = append(current.Hunks, *currentHunk)

		case strings.HasPrefix(line, "+"):
			if current != nil && currentHunk != nil {
				idx := len(current.Hunks) - 1
				current.Hunks[idx].Lines = append(current.Hunks[idx].Lines, DiffLine{
					Kind:    LineInsert,
					Content: line[1:],
					NewNum:  current.Hunks[idx].NewStart,
				})
				current.Hunks[idx].NewStart++
			}

		case strings.HasPrefix(line, "-"):
			if current != nil && currentHunk != nil {
				idx := len(current.Hunks) - 1
				current.Hunks[idx].Lines = append(current.Hunks[idx].Lines, DiffLine{
					Kind:    LineDelete,
					Content: line[1:],
					OldNum:  current.Hunks[idx].OldStart,
				})
				current.Hunks[idx].OldStart++
			}

		default:
			// Context line (starts with space) or blank line
			content := line
			if strings.HasPrefix(line, " ") {
				content = line[1:]
			}
			if current != nil && currentHunk != nil {
				// Skip blank lines that aren't part of the diff content
				if strings.TrimSpace(content) == "" && strings.TrimSpace(line) == "" {
					continue
				}
				idx := len(current.Hunks) - 1
				current.Hunks[idx].Lines = append(current.Hunks[idx].Lines, DiffLine{
					Kind:    LineContext,
					Content: content,
					OldNum:  current.Hunks[idx].OldStart,
					NewNum:  current.Hunks[idx].NewStart,
				})
				current.Hunks[idx].OldStart++
				current.Hunks[idx].NewStart++
			}
		}
	}

	if current != nil {
		files = append(files, *current)
	}
	return files
}

func parseHunkHeader(line string) (int, int) {
	// Parse "@@ -oldStart,oldCount +newStart,newCount @@"
	oldStart, newStart := 1, 1
	line = strings.TrimPrefix(line, "@@ ")
	if idx := strings.Index(line, " @@"); idx >= 0 {
		line = line[:idx]
	}
	parts := strings.Fields(line)
	for _, p := range parts {
		p = strings.TrimPrefix(p, "-")
		p = strings.TrimPrefix(p, "+")
		if idx := strings.IndexByte(p, ','); idx >= 0 {
			p = p[:idx]
		}
		if n := atoi(p); n > 0 {
			if strings.Contains(p, "-") || oldStart == 1 {
				// heuristic: first number is oldStart
			}
		}
	}
	// Simpler approach: extract numbers
	for i, p := range parts {
		clean := strings.TrimLeft(p, "-+")
		if idx := strings.IndexByte(clean, ','); idx >= 0 {
			clean = clean[:idx]
		}
		n := atoi(clean)
		if i == 0 && n > 0 {
			oldStart = n
		} else if i == 1 && n > 0 {
			newStart = n
		}
	}
	return oldStart, newStart
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}
