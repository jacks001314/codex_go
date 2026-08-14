package tui

import (
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	InlineVisualizationDirectivePrefix = "::codex-inline-vis{"
	maxInlineVisualizationBytes        = 2 * 1024 * 1024
)

var inlineVisualizationDirective = regexp.MustCompile(`^::codex-inline-vis\{file="([^"]+)"\}\s*$`)

type InlineVisualization struct {
	Label string
	URL   string
}

type InlineVisualizationContext struct {
	VisualizationsDir string
	ThreadDir         string
	// ViewerDir is a dedicated cache, outside the visualization artifacts,
	// where browser viewer documents are materialized (Rust #38306). When empty
	// it falls back to the legacy `.codex-viewers` directory beside the
	// artifact thread directory.
	ViewerDir string
	// DisableViewers disables link generation entirely. Callers set this when
	// the active filesystem policy can write to the viewer cache (for example a
	// full-disk-write session), so materialized viewer documents cannot be
	// tampered with before they are opened in a browser (Rust #38306).
	DisableViewers bool
}

// RewriteInlineVisualizations converts committed directives to browser links. Incomplete
// directives are hidden while streaming, and unavailable artifacts get an explicit fallback.
func RewriteInlineVisualizations(markdown string, context *InlineVisualizationContext) (string, []InlineVisualization) {
	if !strings.Contains(markdown, InlineVisualizationDirectivePrefix) {
		return markdown, nil
	}
	lines := strings.SplitAfter(markdown, "\n")
	var out strings.Builder
	visualizations := []InlineVisualization{}
	inFence := false
	for _, sourceLine := range lines {
		line := strings.TrimSuffix(sourceLine, "\n")
		newline := ""
		if strings.HasSuffix(sourceLine, "\n") {
			newline = "\n"
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			out.WriteString(line)
			out.WriteString(newline)
			continue
		}
		if inFence || !strings.HasPrefix(trimmed, InlineVisualizationDirectivePrefix) {
			out.WriteString(line)
			out.WriteString(newline)
			continue
		}
		match := inlineVisualizationDirective.FindStringSubmatch(trimmed)
		if len(match) != 2 {
			if strings.HasSuffix(trimmed, "}") {
				out.WriteString("_Visualization unavailable on this device._")
			}
			out.WriteString(newline)
			continue
		}
		viewerURL, ok := context.viewerURL(match[1])
		if !ok {
			out.WriteString("_Visualization unavailable on this device._")
			out.WriteString(newline)
			continue
		}
		name := strings.TrimSuffix(filepath.Base(match[1]), filepath.Ext(match[1]))
		if name == "" {
			name = "generated"
		}
		label := fmt.Sprintf("Open %s visualization in the browser", name)
		out.WriteString(label)
		out.WriteString("  \n")
		out.WriteString("[")
		out.WriteString(viewerURL)
		out.WriteString("](")
		out.WriteString(viewerURL)
		out.WriteString(")")
		out.WriteString(newline)
		visualizations = append(visualizations, InlineVisualization{Label: label, URL: viewerURL})
	}
	return out.String(), visualizations
}

func ExtractInlineVisualizations(markdown string) (string, []InlineVisualization) {
	return RewriteInlineVisualizations(markdown, nil)
}

func (context *InlineVisualizationContext) viewerURL(file string) (string, bool) {
	if context == nil || filepath.Ext(file) != ".html" || filepath.Base(file) != file {
		return "", false
	}
	if context.DisableViewers {
		return "", false
	}
	visualizationsDir, err := filepath.EvalSymlinks(context.VisualizationsDir)
	if err != nil {
		return "", false
	}
	threadDir, err := filepath.EvalSymlinks(context.ThreadDir)
	if err != nil || !pathWithin(threadDir, visualizationsDir) {
		return "", false
	}
	fragmentPath, err := filepath.EvalSymlinks(filepath.Join(threadDir, file))
	if err != nil || !pathWithin(fragmentPath, threadDir) {
		return "", false
	}
	info, err := os.Stat(fragmentPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxInlineVisualizationBytes {
		return "", false
	}
	fragment, err := os.ReadFile(fragmentPath)
	if err != nil {
		return "", false
	}
	viewerDir := filepath.Join(threadDir, ".codex-viewers")
	if strings.TrimSpace(context.ViewerDir) != "" {
		viewerDir = context.ViewerDir
	}
	if err := os.MkdirAll(viewerDir, 0o700); err != nil {
		return "", false
	}
	viewerPath := filepath.Join(viewerDir, file)
	document := inlineVisualizationViewerDocument(string(fragment), strings.TrimSuffix(file, ".html"))
	if existing, readErr := os.ReadFile(viewerPath); readErr != nil || string(existing) != document {
		temporary, createErr := os.CreateTemp(viewerDir, ".viewer-*")
		if createErr != nil {
			return "", false
		}
		temporaryPath := temporary.Name()
		defer os.Remove(temporaryPath)
		if _, err = temporary.WriteString(document); err == nil {
			err = temporary.Close()
		} else {
			_ = temporary.Close()
		}
		if err != nil || os.Rename(temporaryPath, viewerPath) != nil {
			return "", false
		}
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(viewerPath)}).String(), true
}

func pathWithin(path string, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func inlineVisualizationViewerDocument(fragment string, title string) string {
	frame := `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline' 'unsafe-eval' blob: data: https:; style-src 'unsafe-inline' blob: data: https:; img-src blob: data: https:; font-src blob: data: https:; connect-src blob: data:; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'"><title>` + html.EscapeString(title) + `</title></head><body>` + fragment + `</body></html>`
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="referrer" content="no-referrer"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; frame-src 'self'"><title>` + html.EscapeString(title) + `</title><style>html,body{margin:0}iframe{width:100%;height:100vh;border:0}</style></head><body><iframe sandbox="allow-scripts" referrerpolicy="no-referrer" srcdoc="` + html.EscapeString(frame) + `"></iframe></body></html>`
}
