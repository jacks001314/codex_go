package tea

import "strings"

// clipOwnedSectionsToHeight mirrors Rust #47096's short-terminal order for the
// owned transcript: shared hints clip before queued/activity previews, and the
// composer and status row keep their rows. Transcript rows shrink last, from the
// top, so the latest conversation content stays visible.
func clipOwnedSectionsToHeight(sections []string, height int, footerIndex, previewIndex, hudIndex, workingIndex, composerIndex *int) []string {
	if height <= 0 || len(sections) == 0 {
		return sections
	}
	total := 0
	for _, section := range sections {
		total += sectionHeight(section)
	}
	overflow := total - height
	if overflow <= 0 {
		return sections
	}
	remove := map[int]bool{}
	indexes := []*int{footerIndex, hudIndex, workingIndex, previewIndex}
	for _, index := range indexes {
		if overflow <= 0 {
			break
		}
		if index == nil || *index < 0 || *index >= len(sections) || remove[*index] {
			continue
		}
		remove[*index] = true
		overflow -= sectionHeight(sections[*index])
	}
	out := make([]string, 0, len(sections))
	remapped := make(map[int]int, len(sections))
	for index, section := range sections {
		if !remove[index] {
			remapped[index] = len(out)
			out = append(out, section)
		}
	}
	tracked := []*int{footerIndex, previewIndex, hudIndex, workingIndex, composerIndex}
	for _, index := range tracked {
		if index == nil || *index < 0 {
			continue
		}
		if next, ok := remapped[*index]; ok {
			*index = next
		} else {
			*index = -1
		}
	}
	if overflow > 0 && len(out) > 1 {
		lines := strings.Split(out[1], "\n")
		cut := min(overflow, max(len(lines)-minTranscriptHeight, 0))
		if cut > 0 {
			out[1] = strings.Join(lines[cut:], "\n")
		}
	}
	return out
}
