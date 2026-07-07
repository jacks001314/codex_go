package review

import (
	"fmt"
	"strings"
)

const FallbackMessage = "Reviewer failed to output a response."

type CodeLocation struct {
	AbsoluteFilePath string
	StartLine        int
	EndLine          int
}

type Finding struct {
	Title        string
	Body         string
	CodeLocation CodeLocation
}

type OutputEvent struct {
	OverallExplanation string
	Findings           []Finding
}

func FormatLocation(item *Finding) string {
	if item == nil {
		return ""
	}
	end := item.CodeLocation.EndLine
	if end == 0 {
		end = item.CodeLocation.StartLine
	}
	return fmt.Sprintf("%s:%d-%d", item.CodeLocation.AbsoluteFilePath, item.CodeLocation.StartLine, end)
}

func FormatFindingsBlock(findings []Finding, selection []bool) string {
	lines := []string{""}
	if len(findings) > 1 {
		lines = append(lines, "Full review comments:")
	} else {
		lines = append(lines, "Review comment:")
	}
	for index := range findings {
		item := &findings[index]
		lines = append(lines, "")
		prefix := "-"
		if selection != nil {
			checked := true
			if index < len(selection) {
				checked = selection[index]
			}
			if checked {
				prefix = "- [x]"
			} else {
				prefix = "- [ ]"
			}
		}
		lines = append(lines, fmt.Sprintf("%s %s - %s", prefix, item.Title, FormatLocation(item)))
		for _, line := range strings.Split(item.Body, "\n") {
			if line == "" {
				lines = append(lines, "  ")
			} else {
				lines = append(lines, "  "+line)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func RenderOutputText(output *OutputEvent) string {
	if output == nil {
		return FallbackMessage
	}
	var sections []string
	if explanation := strings.TrimSpace(output.OverallExplanation); explanation != "" {
		sections = append(sections, explanation)
	}
	if len(output.Findings) > 0 {
		sections = append(sections, strings.TrimSpace(FormatFindingsBlock(output.Findings, nil)))
	}
	if len(sections) == 0 {
		return FallbackMessage
	}
	return strings.Join(sections, "\n\n")
}
