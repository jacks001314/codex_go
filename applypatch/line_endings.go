package applypatch

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
)

// PreserveLineEndingsEnvVar carries the selected apply_patch update mode
// through arg0-dispatched standalone apply_patch processes. Mirrors Rust
// CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS_ENV_VAR (21aa552e87).
const PreserveLineEndingsEnvVar = "CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS"

// FileUpdateMode controls how updates reconstruct the target file after
// matching a patch. Mirrors Rust ApplyPatchFileUpdateMode (21aa552e87).
type FileUpdateMode int

const (
	// UpdateModeNormalizeToLF preserves the historical behavior of
	// normalizing updated files to LF.
	UpdateModeNormalizeToLF FileUpdateMode = iota
	// UpdateModePreserveLineEndings preserves existing line endings and uses
	// the file's preferred ending for new lines.
	UpdateModePreserveLineEndings
)

// FileUpdateModeFromEnv reads the update mode selected for an
// arg0-dispatched apply_patch process.
func FileUpdateModeFromEnv() FileUpdateMode {
	if os.Getenv(PreserveLineEndingsEnvVar) == "1" {
		return UpdateModePreserveLineEndings
	}
	return UpdateModeNormalizeToLF
}

type lineEnding int

const (
	lineEndingLF lineEnding = iota
	lineEndingCRLF
	lineEndingCR
)

func (e lineEnding) String() string {
	switch e {
	case lineEndingCRLF:
		return "\r\n"
	case lineEndingCR:
		return "\r"
	default:
		return "\n"
	}
}

type sourceLine struct {
	text   string
	ending *lineEnding
}

// sourceFile mirrors Rust text_file::SourceFile (21aa552e87): a logical view
// of the file contents that retains each line's ending.
type sourceFile struct {
	lines           []sourceLine
	preferredEnding lineEnding
}

// parseSourceFile splits contents into logical lines while retaining each
// line ending. The first existing ending becomes the preferred style for
// inserted lines; files without an ending default to LF.
func parseSourceFile(contents string) *sourceFile {
	var lines []sourceLine
	var preferred *lineEnding
	lineStart := 0
	cursor := 0
	for cursor < len(contents) {
		var ending lineEnding
		var endingLen int
		switch {
		case contents[cursor] == '\r' && cursor+1 < len(contents) && contents[cursor+1] == '\n':
			ending, endingLen = lineEndingCRLF, 2
		case contents[cursor] == '\r':
			ending, endingLen = lineEndingCR, 1
		case contents[cursor] == '\n':
			ending, endingLen = lineEndingLF, 1
		default:
			cursor++
			continue
		}
		if preferred == nil {
			value := ending
			preferred = &value
		}
		lines = append(lines, sourceLine{text: contents[lineStart:cursor], ending: &ending})
		cursor += endingLen
		lineStart = cursor
	}
	if lineStart < len(contents) {
		lines = append(lines, sourceLine{text: contents[lineStart:], ending: nil})
	}
	preferredEnding := lineEndingLF
	if preferred != nil {
		preferredEnding = *preferred
	}
	return &sourceFile{lines: lines, preferredEnding: preferredEnding}
}

func (f *sourceFile) lineTexts() []string {
	texts := make([]string, 0, len(f.lines))
	for _, line := range f.lines {
		texts = append(texts, line.text)
	}
	return texts
}

type replacement struct {
	start    int
	oldLen   int
	newLines []string
}

// applyReplacements rebuilds the file from source-ordered, non-overlapping
// replacements. Unchanged lines retain their original endings, inserted lines
// use the preferred ending, and every resulting line receives an ending to
// match apply-patch's historical trailing-newline behavior.
func (f *sourceFile) applyReplacements(replacements []replacement) {
	original := f.lines
	var newLines []sourceLine
	sourceIndex := 0
	for _, rep := range replacements {
		if rep.start < sourceIndex {
			continue
		}
		newLines = append(newLines, original[sourceIndex:rep.start]...)
		for _, text := range rep.newLines {
			ending := f.preferredEnding
			newLines = append(newLines, sourceLine{text: text, ending: &ending})
		}
		sourceIndex = rep.start + rep.oldLen
	}
	newLines = append(newLines, original[sourceIndex:]...)
	f.lines = newLines

	// Updates have historically added a trailing newline. This also gives an
	// unterminated last line an ending if an insertion moved it inward.
	for i := range f.lines {
		if f.lines[i].ending == nil {
			ending := f.preferredEnding
			f.lines[i].ending = &ending
		}
	}
}

func (f *sourceFile) intoContents() string {
	var builder strings.Builder
	for _, line := range f.lines {
		builder.WriteString(line.text)
		if line.ending != nil {
			builder.WriteString(line.ending.String())
		}
	}
	return builder.String()
}

// seekSequence mirrors Rust seek_sequence (21aa552e87): find pattern lines
// within lines beginning at or after start, trying exact match, then ignoring
// trailing whitespace, then leading and trailing whitespace, then normalized
// Unicode punctuation. When eof is true the search anchors at the end of the
// file first (preserve mode keeps the start bound).
func seekSequence(lines []string, pattern []string, start int, eof bool, preserve bool) int {
	if len(pattern) == 0 {
		return start
	}
	if len(pattern) > len(lines) {
		return -1
	}
	searchStart := start
	if eof && len(lines) >= len(pattern) {
		eofStart := len(lines) - len(pattern)
		if preserve {
			if eofStart > start {
				searchStart = eofStart
			}
		} else {
			searchStart = eofStart
		}
	}
	limit := len(lines) - len(pattern)

	for i := searchStart; i <= limit; i++ {
		match := true
		for j := range pattern {
			if lines[i+j] != pattern[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	for i := searchStart; i <= limit; i++ {
		match := true
		for j := range pattern {
			if strings.TrimRightFunc(lines[i+j], unicode.IsSpace) != strings.TrimRightFunc(pattern[j], unicode.IsSpace) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	for i := searchStart; i <= limit; i++ {
		match := true
		for j := range pattern {
			if strings.TrimSpace(lines[i+j]) != strings.TrimSpace(pattern[j]) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	for i := searchStart; i <= limit; i++ {
		match := true
		for j := range pattern {
			if normalizeSearchLine(lines[i+j]) != normalizeSearchLine(pattern[j]) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// normalizeSearchLine mirrors Rust seek_sequence::normalise: trim and map
// typographic punctuation to ASCII equivalents so diffs authored with plain
// ASCII can still apply to files with fancy dashes / quotes.
func normalizeSearchLine(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range strings.TrimSpace(value) {
		switch r {
		case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
			builder.WriteRune('-')
		case '\u2018', '\u2019', '\u201A', '\u201B':
			builder.WriteRune('\'')
		case '\u201C', '\u201D', '\u201E', '\u201F':
			builder.WriteRune('"')
		case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007',
			'\u2008', '\u2009', '\u200A', '\u202F', '\u205F', '\u3000':
			builder.WriteRune(' ')
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// applyUnifiedDiffToContentPreserving applies the patch while retaining the
// original line endings of untouched and context lines, using the file's first
// line ending for inserted or replaced lines.
func applyUnifiedDiffToContentPreserving(content string, diff string) (string, error) {
	chunks, err := parseUpdateChunks(diff)
	if err != nil {
		return "", err
	}
	if len(chunks) == 0 {
		return "", fmt.Errorf("%w: empty update hunk", ErrInvalidPatch)
	}
	file := parseSourceFile(content)
	originalLines := file.lineTexts()
	replacements, err := computeReplacementsPreserving(originalLines, chunks)
	if err != nil {
		return "", err
	}
	file.applyReplacements(replacements)
	return file.intoContents(), nil
}

// computeReplacementsPreserving mirrors Rust file_update::compute_replacements
// in PreserveLineEndings mode: context lines occur on both sides of a patch
// chunk and are kept in place so their exact contents and terminators survive,
// especially in mixed-ending files.
func computeReplacementsPreserving(originalLines []string, chunks []*updateChunk) ([]replacement, error) {
	var replacements []replacement
	lineIndex := 0
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		if strings.TrimSpace(chunk.changeContext) != "" {
			idx := seekSequence(originalLines, []string{chunk.changeContext}, lineIndex, false, true)
			if idx < 0 {
				return nil, fmt.Errorf("failed to find context '%s'", chunk.changeContext)
			}
			lineIndex = idx + 1
		}
		if len(chunk.oldLines) == 0 {
			replacements = append(replacements, replacement{
				start:    len(originalLines),
				oldLen:   0,
				newLines: append([]string(nil), chunk.newLines...),
			})
			continue
		}

		pattern := chunk.oldLines
		newSlice := chunk.newLines
		found := seekSequence(originalLines, pattern, lineIndex, chunk.isEndOfFile, true)
		if found < 0 && len(pattern) > 0 && pattern[len(pattern)-1] == "" {
			// Retry without the trailing empty line that represents the final
			// newline in the file.
			pattern = pattern[:len(pattern)-1]
			if len(newSlice) > 0 && newSlice[len(newSlice)-1] == "" {
				newSlice = newSlice[:len(newSlice)-1]
			}
			found = seekSequence(originalLines, pattern, lineIndex, chunk.isEndOfFile, true)
		}
		if found < 0 {
			return nil, fmt.Errorf("failed to find expected lines:\n%s", strings.Join(chunk.oldLines, "\n"))
		}

		oldStart := 0
		newStart := 0
		for _, pair := range chunk.contextIndices {
			oldCtx, newCtx := pair[0], pair[1]
			// A trailing empty context line can be removed from pattern and
			// newSlice above when it represents the final newline.
			if oldCtx >= len(pattern) || newCtx >= len(newSlice) {
				break
			}
			if oldStart != oldCtx || newStart != newCtx {
				replacements = append(replacements, replacement{
					start:    found + oldStart,
					oldLen:   oldCtx - oldStart,
					newLines: append([]string(nil), newSlice[newStart:newCtx]...),
				})
			}
			oldStart = oldCtx + 1
			newStart = newCtx + 1
		}
		if oldStart != len(pattern) || newStart != len(newSlice) {
			replacements = append(replacements, replacement{
				start:    found + oldStart,
				oldLen:   len(pattern) - oldStart,
				newLines: append([]string(nil), newSlice[newStart:]...),
			})
		}
		lineIndex = found + len(pattern)
	}
	sort.SliceStable(replacements, func(i, j int) bool {
		return replacements[i].start < replacements[j].start
	})
	return replacements, nil
}
