package historycell

import (
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/patches.rs.

type PatchHistoryCell struct {
	Changes map[string]tui.FileChange
	CWD     string
	Theme   string
}

func NewPatchEvent(changes map[string]tui.FileChange, cwd string) PatchHistoryCell {
	return newPatchEventTheme(changes, cwd, "dark")
}

func NewPatchEventWithTheme(changes map[string]tui.FileChange, cwd string, theme string) PatchHistoryCell {
	return newPatchEventTheme(changes, cwd, theme)
}

func newPatchEventTheme(changes map[string]tui.FileChange, cwd string, theme string) PatchHistoryCell {
	cloned := make(map[string]tui.FileChange, len(changes))
	for path, change := range changes {
		cloned[path] = change
	}
	return PatchHistoryCell{Changes: cloned, CWD: strings.TrimSpace(cwd), Theme: theme}
}

func (c PatchHistoryCell) DisplayLines(width int) []string {
	// Rust #42107: TUI history renders the complete patch inline.
	return tui.CreateDiffSummary(c.Changes, c.CWD, width, c.Theme)
}

func (c PatchHistoryCell) RawLines() []string {
	return tui.CreateDiffSummary(c.Changes, c.CWD, 120, c.Theme)
}

func NewPatchApplyFailure(stderr string) PlainHistoryCell {
	return NewPlainHistoryCell(patchApplyFailureLines(stderr, 5))
}

func patchApplyFailureLines(stderr string, lineLimit int) []string {
	lines := strings.Split(strings.ReplaceAll(stderr, "\r\n", "\n"), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if lineLimit < 1 {
		lineLimit = 1
	}
	selected := lines
	if len(lines) > lineLimit*2 {
		omitted := len(lines) - lineLimit*2
		selected = append([]string(nil), lines[:lineLimit]...)
		label := "… +" + strconv.Itoa(omitted) + " lines"
		if hint := tui.TranscriptDisclosureHint(); hint != "" {
			label += " (" + hint + ")"
		}
		selected = append(selected, label)
		selected = append(selected, lines[len(lines)-lineLimit:]...)
	}
	out := []string{"\u2718 Failed to apply patch"}
	for _, line := range selected {
		prefix := "  "
		if len(out) == 1 {
			prefix = "| "
		}
		out = append(out, prefix+line)
	}
	return out
}

// ViewImageHistoryCell renders a viewed local image and carries the artifact
// path the owned fullscreen transcript may preview.
type ViewImageHistoryCell struct {
	ActivityID string
	Path       string
	Label      string
	CWD        string
	// LinkDestination is the OSC-8 file destination of the displayed image path
	// (Rust ViewImageHistoryCell::link, #58446). Empty means the row is plain
	// text: the workspace is remote, the session is remote, or the path is not a
	// local regular image file.
	LinkDestination string
}

// LinkLocalImage attaches the file hyperlink for a locally viewed image when the
// path resolves to a regular image file (Rust link_local_image). The caller
// decides whether the workspace is local and the session is not an SSH one.
func (c ViewImageHistoryCell) LinkLocalImage() ViewImageHistoryCell {
	if destination, ok := tui.ViewedImageDestination(c.Path); ok {
		c.LinkDestination = destination
	}
	return c
}

// NewViewImageToolCall creates a viewed-image cell for callers without an item
// id. The path-derived identity keeps the preview behind disclosure, matching
// Rust's always-identified ImageView cell.
func NewViewImageToolCall(path string, cwd string) ViewImageHistoryCell {
	return NewViewImageToolCallWithID("legacy:"+strings.TrimSpace(path), path, cwd)
}

// NewViewImageToolCallWithID creates a viewed-image cell whose activity ID
// controls disclosure of the optional preview.
func NewViewImageToolCallWithID(id string, path string, cwd string) ViewImageHistoryCell {
	cwd = strings.TrimSpace(cwd)
	return ViewImageHistoryCell{
		ActivityID: strings.TrimSpace(id),
		Path:       absoluteImagePath(path, cwd),
		Label:      strings.TrimSpace(path),
		CWD:        cwd,
	}
}

// displayFilename is the compact row's file name: the path relative to the
// session cwd when it stays inside it, otherwise the label as given.
func (c ViewImageHistoryCell) displayFilename() string {
	displayPath := strings.TrimSpace(c.Label)
	if displayPath != "" && c.CWD != "" {
		if abs, err := filepath.Abs(c.Path); err == nil {
			if rel, err := filepath.Rel(c.CWD, abs); err == nil && !strings.HasPrefix(rel, "..") {
				displayPath = filepath.ToSlash(rel)
			}
		}
	}
	if displayPath == "" {
		displayPath = c.Label
	}
	filename := strings.TrimRight(displayPath, `/\`)
	if index := strings.LastIndexAny(filename, `/\`); index >= 0 {
		filename = filename[index+1:]
	}
	if filename == "" {
		filename = displayPath
	}
	return strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(filename)
}

// DisplayHyperlinkLines renders the compact row and keeps the image path linked
// through truncation (Rust display_hyperlink_lines, #58446).
func (c ViewImageHistoryCell) DisplayHyperlinkLines(width int) []HyperlinkLine {
	line := HyperlinkLine{Text: "\u2022 Viewed image " + c.displayFilename()}
	line.Links = c.linkWithin(line.Text, tui.DisplayWidth("\u2022 Viewed image "))
	if width > 0 {
		line = ClipHyperlinkLine(line, width)
	}
	return []HyperlinkLine{line}
}

func (c ViewImageHistoryCell) DisplayLines(width int) []string {
	return RenderHyperlinkLines(c.DisplayHyperlinkLines(width))
}

func (c ViewImageHistoryCell) RawLines() []string {
	path := strings.TrimSpace(c.Label)
	if path == "" {
		path = c.Path
	}
	return []string{"Viewed image " + path}
}

// transcriptPath is the complete image path shown in the expanded transcript.
func (c ViewImageHistoryCell) transcriptPath() string {
	path := strings.TrimSpace(c.Label)
	if path == "" {
		path = c.Path
	}
	return path
}

// linkWithin returns the image link over the path portion of a rendered row.
func (c ViewImageHistoryCell) linkWithin(text string, start int) []tui.TerminalHyperlink {
	if strings.TrimSpace(c.LinkDestination) == "" {
		return nil
	}
	if end := tui.DisplayWidth(text); end > start {
		return []tui.TerminalHyperlink{{Start: start, End: end, Destination: c.LinkDestination}}
	}
	return nil
}

// TranscriptHyperlinkLines keeps the complete image path linked while the
// transcript wraps it (Rust transcript_hyperlink_lines, #58446).
func (c ViewImageHistoryCell) TranscriptHyperlinkLines(width int) []HyperlinkLine {
	line := HyperlinkLine{Text: "Viewed image " + c.transcriptPath()}
	line.Links = c.linkWithin(line.Text, tui.DisplayWidth("Viewed image "))
	if width <= 0 {
		return []HyperlinkLine{line}
	}
	return AdaptiveWrapHyperlinkLines([]HyperlinkLine{line}, tui.WrapOptions{
		Width:            width,
		InitialIndent:    "\u2022 ",
		SubsequentIndent: "  ",
		BreakWords:       true,
	})
}

// TranscriptLines retains the complete image path in the expanded transcript
// (Rust #46116 transcript_lines).
func (c ViewImageHistoryCell) TranscriptLines(width int) []string {
	return RenderHyperlinkLines(c.TranscriptHyperlinkLines(width))
}

// ActivityIDs returns the stable disclosure identity for the viewed image.
func (c ViewImageHistoryCell) ActivityIDs() []string {
	if c.ActivityID == "" {
		return nil
	}
	return []string{"image-view:" + c.ActivityID}
}

// ActivityDisclosure reports generic transcript details for a viewed image
// (Rust's default HistoryCell::activity_disclosure).
func (c ViewImageHistoryCell) ActivityDisclosure(int) tui.ActivityDisclosure {
	return tui.ActivityDisclosure{Generic: true}
}

// ImagePath returns the local artifact path for transcript previews.
func (c ViewImageHistoryCell) ImagePath() string {
	return c.Path
}

// ImageGenerationHistoryCell renders a generated-image result and carries the
// saved local artifact for transcript previews.
type ImageGenerationHistoryCell struct {
	Lines     []string
	SavedPath string
}

// NewImageGenerationCall creates a generated-image cell. Failed generations
// keep the saved-path text but do not offer a preview.
func NewImageGenerationCall(callID string, status string, revisedPrompt string, savedPath string) ImageGenerationHistoryCell {
	detail := firstNonEmptyHistory(revisedPrompt, callID)
	heading := "\u2022 Generated Image:"
	failed := strings.EqualFold(strings.TrimSpace(status), "failed")
	if failed {
		heading = "\u2717 Image generation failed"
	}
	lines := []string{heading, "  \u2514 " + detail}
	previewPath := ""
	if strings.TrimSpace(savedPath) != "" {
		lines = append(lines, "  \u2514 Saved to: "+strings.TrimSpace(savedPath))
		if !failed {
			previewPath = absoluteImagePath(savedPath, "")
		}
	}
	return ImageGenerationHistoryCell{Lines: lines, SavedPath: previewPath}
}

func (c ImageGenerationHistoryCell) DisplayLines(width int) []string {
	_ = width
	return append([]string(nil), c.Lines...)
}

func (c ImageGenerationHistoryCell) RawLines() []string {
	return plainLines(c.Lines)
}

// ImagePath returns the saved local artifact path for transcript previews.
func (c ImageGenerationHistoryCell) ImagePath() string {
	return c.SavedPath
}

// absoluteImagePath resolves a local artifact (including a file:// URL) against
// the supplied cwd so decoding never depends on the process working directory.
func absoluteImagePath(path string, cwd string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if parsed, err := url.Parse(path); err == nil && parsed.Scheme == "file" {
		path = parsed.Path
		if parsed.Host != "" && parsed.Host != "localhost" {
			path = "//" + parsed.Host + parsed.Path
		}
		if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
			path = path[1:]
		}
		path = filepath.FromSlash(path)
	}
	if !filepath.IsAbs(path) {
		if cwd == "" {
			if abs, err := filepath.Abs(path); err == nil {
				path = abs
			}
		} else {
			path = filepath.Join(cwd, path)
		}
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
