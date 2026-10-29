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

// clipSectionsToHeight mirrors Rust's flex layout for the owned screen: the
// transcript yields rows first, then the flexible activity/preview rows, and
// only then does the composer lose rows. Sections removed here are unlinked
// from the tracked indices so later selection registration stays accurate.
func clipSectionsToHeight(
	sections []string,
	height int,
	transcriptIndex *int,
	composerIndex *int,
	statusIndex *int,
	footerIndex *int,
	workingIndex *int,
	hudIndex *int,
	previewIndex *int,
	warningBodyIndex *int,
) []string {
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
	store := map[int]string{}
	valid := func(index *int) bool {
		return index != nil && *index >= 0 && *index < len(sections) && !remove[*index]
	}
	if valid(transcriptIndex) {
		lines := strings.Split(sections[*transcriptIndex], "\n")
		if len(lines) <= overflow {
			remove[*transcriptIndex] = true
			overflow -= len(lines)
		} else {
			store[*transcriptIndex] = strings.Join(lines[overflow:], "\n")
			overflow = 0
		}
	}
	for _, index := range []*int{workingIndex, hudIndex, previewIndex} {
		if overflow <= 0 {
			break
		}
		if !valid(index) {
			continue
		}
		remove[*index] = true
		overflow -= sectionHeight(sections[*index])
	}
	// The blank gap row above the composer is the next flexible row.
	if overflow > 0 && composerIndex != nil {
		for index := *composerIndex - 1; index >= 0; index-- {
			if remove[index] || strings.TrimSpace(sections[index]) != "" {
				continue
			}
			remove[index] = true
			overflow -= 1
			break
		}
	}
	if overflow > 0 && valid(composerIndex) {
		lines := strings.Split(sections[*composerIndex], "\n")
		cut := min(overflow, max(len(lines)-1, 0))
		if cut > 0 {
			store[*composerIndex] = strings.Join(lines[:len(lines)-cut], "\n")
			overflow -= cut
		}
	}
	out := make([]string, 0, len(sections))
	remapped := make(map[int]int, len(sections))
	for index, section := range sections {
		if remove[index] {
			continue
		}
		if replacement, ok := store[index]; ok {
			section = replacement
		}
		remapped[index] = len(out)
		out = append(out, section)
	}
	for _, index := range []*int{transcriptIndex, composerIndex, statusIndex, footerIndex, workingIndex, hudIndex, previewIndex, warningBodyIndex} {
		if index == nil || *index < 0 {
			continue
		}
		if next, ok := remapped[*index]; ok {
			*index = next
		} else {
			*index = -1
		}
	}
	return out
}
